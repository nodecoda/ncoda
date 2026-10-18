//@feature hoist-zero-default — 反编译提升变量补类型零值（dify runtime 缺省值安全网显式化，DIFY-DECOMPILE §0）

package decompile

// round2_regression_test.go —— 分支处理回归测试（2026-08-29 继承 coze-biz
// 分支处理经验后的定向锁定）:
//   1. 提升变量 declared 基集注册 → answer 引用分支内节点可解析（03）;
//   2. 未引用分支产值 → 裸 ExprStmt（03 的 false 分支 llm 不声明变量）;
//   3. barrier 组合 → 分支走到外层收敛点即停,不重复转换共享终点
//      （test_complex_branch 的 parallel 兄弟分支读写竞争）。
// 断言反编译产物语义形态;compile_ok 由 TestRound2FixedPoint 全量门禁覆盖。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decompileFixture(t *testing.T, name string) string {
	t.Helper()
	dir := os.Getenv("DIFY_YAML_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "examples", "yaml")
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("%s: read: %v", name, err)
	}
	nc, err := DecompileYAML(string(b))
	if err != nil {
		t.Fatalf("%s: decompile: %v", name, err)
	}
	return nc
}

// TestRound2HoistedVarResolved 提升变量注册进 declared 基集后,分支之后的
// answer 引用必须解析到提升变量,而不是丢失为 return ""。
func TestRound2HoistedVarResolved(t *testing.T) {
	nc := decompileFixture(t, "03_conditional_branch.yml")
	if !strings.Contains(nc, "return vllm_zh.text;") {
		t.Errorf("answer 引用 llm-zh 应解析为 return vllm_zh.text;,实际:\n%s", nc)
	}
	if strings.Contains(nc, "return \"\";") {
		t.Errorf("answer 引用不得丢失为 return \"\";实际:\n%s", nc)
	}
}

// TestRound2UnusedBranchVarDiscarded 产值节点若未被任何下游引用 → 丢弃为
// 裸表达式语句（不声明变量）,否则 UNUSED_VAR 升级 ANALYSIS_ERROR。
// 99_dead_nodes 的 llm 无任何下游引用 → 裸调用 llm(MODEL_1, {}) + return ""。
func TestRound2UnusedBranchVarDiscarded(t *testing.T) {
	nc := decompileFixture(t, "99_dead_nodes.yml")
	if strings.Contains(nc, "let v") {
		t.Errorf("未引用的产值节点不得声明变量,应裸调用;实际:\n%s", nc)
	}
	// 裸调用保留副作用:仍有 llm(MODEL_1, {}) 调用。
	if !strings.Contains(nc, "llm(MODEL_1,") {
		t.Errorf("未引用 llm 调用应保留（副作用）;实际:\n%s", nc)
	}
}

// TestRound2AnswerTerminalRefDeclaresBranchVar 03 的 else 分支 llm 输出虽
// 未被 answer 模板字面引用,但 answer 在该路径经 pathTerminal 重定向到本
// 路径末端 producer（vllm_en）→ 必须声明变量,否则 return vllm_en.text
// 引用未声明变量 → UNDEFINED_VAR。这是 collectTerminalRefs 的回归锚点
// （collectRefs 漏掉 pathTerminal 隐式引用 → 误判未引用 → 降级裸调用）。
func TestRound2AnswerTerminalRefDeclaresBranchVar(t *testing.T) {
	nc := decompileFixture(t, "03_conditional_branch.yml")
	// else 分支声明 vllm_en 并 return vllm_en.text（answer 经 pathTerminal
	// 引用本路径末端 producer——true 分支引用 llm-zh,false 分支引用 llm-en）。
	if !strings.Contains(nc, "let vllm_en = llm(MODEL_2,") {
		t.Errorf("else 分支 llm 输出被 answer 经 pathTerminal 引用,必须声明 vllm_en;实际:\n%s", nc)
	}
	if !strings.Contains(nc, "return vllm_en.text;") {
		t.Errorf("else 分支 answer 引用应解析为 return vllm_en.text;实际:\n%s", nc)
	}
	// true 分支对应 vllm_zh（answer 模板字面引用 llm-zh）。
	if !strings.Contains(nc, "return vllm_zh.text;") {
		t.Errorf("true 分支 answer 引用应解析为 return vllm_zh.text;实际:\n%s", nc)
	}
}

// TestRound2ParallelBarrierNoSiblingHazard 内层 if-else conv 为空时保留
// 外层 barrier:分支走到外层收敛点（answer）即停,不重复转换——否则 parallel
// 分支内引用兄弟分支提升变量 → P5 读写竞争 E1036。
func TestRound2ParallelBarrierNoSiblingHazard(t *testing.T) {
	nc := decompileFixture(t, "test_complex_branch.yml")
	// 分支 1 的 if-else 只保留 true 分支（return "contains 'hello'"）;
	// false 分支在 parallel 内不得出现 return v1755502777322.text。
	pIdx := strings.Index(nc, "parallel {")
	eIdx := strings.Index(nc[pIdx:], "}")
	if pIdx < 0 {
		t.Fatalf("产物缺少 parallel;实际:\n%s", nc)
	}
	inner := nc[pIdx : pIdx+eIdx]
	if strings.Contains(inner, "v1755502777322.text") {
		t.Errorf("parallel 内分支不得引用兄弟分支提升变量（P5 竞争）;parallel 块:\n%s", inner)
	}
	// 收敛点 answer 在 parallel 之后只转一次。
	if !strings.Contains(nc, "return v1755502777322.text;") {
		t.Errorf("parallel 之后应有一次 answer 引用 v1755502777322.text;实际:\n%s", nc)
	}
}

// TestRound2HoistedVarZeroValueInit conditional_parallel 提升变量补类型零值
// 初始（dify runtime 缺省值安全网显式化,见 docs/DIFY-DECOMPILE.md §0）:
//  1. 提升变量带类型声明（Hoist<id> v<id>;）且紧跟 v<id> = <零值>; 赋值
//     —— definite-init 视为已初始化（否则 UNINITIALIZED_USE）;
//  2. 零值形状 = record 全字段零值（string → ""）;
//  3. 分支内赋值覆盖零值,return dict 引用全部可解析。
//
// 语义对齐 dify runtime:end 引用未执行节点输出 = null（graphon 兜底）,
// 反编译产物用类型零值显式化该安全网。
func TestRound2HoistedVarZeroValueInit(t *testing.T) {
	nc := decompileFixture(t, "conditional_parallel_code_execution_workflow.yml")
	// 三个提升变量（code1/code2/code3）都必须带零值初始赋值。
	for _, id := range []string{"v1754230738434", "v17542307611100", "v17542307643480"} {
		if !strings.Contains(nc, id+" = { \"result\": \"\" };") {
			t.Errorf("提升变量 %s 缺少类型零值初始（应为 %s = { \"result\": \"\" };）;产物:\n%s", id, id, nc)
		}
		// 带类型声明必须在零值赋值之前出现（Hoist<id> v<id>;）。
		td := "Hoist" + strings.TrimPrefix(id, "v")
		if !strings.Contains(nc, td+" "+id+";") {
			t.Errorf("提升变量 %s 缺少带类型声明 %s %s;;产物:\n%s", id, td, id, nc)
		}
	}
	// return dict 引用三个提升变量,全部初始化后可解析。
	if !strings.Contains(nc, "return { \"1\": v1754230738434.result, \"2\": v17542307611100.result, \"3\": v17542307643480.result };") {
		t.Errorf("return dict 引用三个提升变量缺失;产物:\n%s", nc)
	}
}
