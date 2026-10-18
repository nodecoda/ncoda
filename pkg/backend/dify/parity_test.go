//go:build shelved

// 搁置面（AGENTS.md「开发主线与平台优先级」2026-09-11 拍板）：dify / coze-studio /
// python golden 语料面的测试结果**不是**主线（coze-biz）回归判据。本文件按门禁治理
// `内部治理文档（G-B 条）` G-B「快照/搁置面不阻塞默认门禁」移到默认门禁之外，
// 以免"常驻红基线"淹没主线真信号（实测：30 条常驻红里 23 条是搁置面）。
//
// 跑它：`go test -tags shelved ./...`（或 `make shelved`）；分类：`scripts/gov/triage.py`。
// 摘掉本 tag 的条件 = 该面重新成为主线判据（届时先修红再摘）。

package dify_test

//@feature attempt

// Golden parity harness: for every corpus file, compare the production
// IR-render output against the frozen legacy-lowering snapshot
// (testdata/parity-golden/<name>.yml). The legacy path was retired
// (2026-08-20): the golden files were generated from the legacy lowering
// output, which the pre-retirement parity harness proved byte-identical to
// the IR render. Any IR lowering regression now turns RED here before the
// platform gates run. Set IR_PARITY_DUMP=1 to write per-case YAMLs to
// /tmp/parity for diffing.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
	"github.com/nodecoda/ncoda/pkg/ir"
	"github.com/nodecoda/ncoda/pkg/passes"
	"github.com/nodecoda/ncoda/pkg/tool"
)

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "parity-golden", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(data)
}

func TestIRRenderParity(t *testing.T) {
	corpus := "../../compiler/testdata/examples/nodecoda"
	files, err := filepath.Glob(filepath.Join(corpus, "*.ncoda"))
	if err != nil {
		t.Fatalf("glob corpus: %v", err)
	}
	// 06_tool_use.ncoda and 38_chatflow_time_tool.ncoda moved to the
	// closed-world qualified-call form in Task 4 (import "dify.google_search";
	// / import "dify.time"; + capability headers). The legacy lowering oracle
	// only dispatches the ident builtins (translate.go dispatch on
	// operation.Name), so no golden snapshot exists for them; they are
	// verified via the dify/coze platform gates and the compiler golden
	// tests instead.
	skipClosedWorld := map[string]bool{
		"06_tool_use.ncoda":           true,
		"38_chatflow_time_tool.ncoda": true,
	}
	dump := os.Getenv("IR_PARITY_DUMP") != ""
	var nPass, nGap, nMismatch, nErr, nSkip int
	for _, f := range files {
		name := filepath.Base(f)
		if skipClosedWorld[name] {
			nSkip++
			t.Logf("SKIP %-42s closed-world qualified-call form (Task 4)", name)
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		res := passes.DefaultPipeline().Run(string(src), name, nil)
		if res.HasErrors() || res.Program == nil {
			nGap++
			t.Logf("GAP  %-42s frontend errors", name)
			continue
		}
		prog := res.Program
		irg, ierr := ir.Build(prog)
		if ierr != nil {
			nGap++
			t.Logf("GAP  %-42s ir-build: %v", name, ierr)
			continue
		}
		gotAST, rerr := dify.RenderFromIR(irg, prog.Program.Mode())
		if rerr != nil {
			nErr++
			t.Logf("ERR  %-42s render: %v", name, rerr)
			continue
		}
		gotYAML := serialize(t, gotAST)
		wantYAML := readGolden(t, name+".yml")
		if gotYAML == wantYAML {
			nPass++
			t.Logf("PASS %-42s", name)
			continue
		}
		nMismatch++
		if dump {
			_ = os.MkdirAll("/tmp/parity", 0o755)
			_ = os.WriteFile("/tmp/parity/"+name+".ir.yml", []byte(gotYAML), 0o644)
			_ = os.WriteFile("/tmp/parity/"+name+".golden.yml", []byte(wantYAML), 0o644)
		}
		t.Logf("DIFF %-42s (IR_PARITY_DUMP=1 → /tmp/parity/%s.*.yml)", name, name)
	}
	t.Logf("=== PASS=%d GAP=%d MISMATCH=%d ERR=%d SKIP=%d (corpus=%d)", nPass, nGap, nMismatch, nErr, nSkip, len(files))
	if nPass+nSkip != len(files) || nGap+nMismatch+nErr > 0 {
		t.Errorf("parity not reached: %d pass, %d gaps, %d mismatches, %d errors, %d skipped", nPass, nGap, nMismatch, nErr, nSkip)
	}
}

func TestIRRenderLoopParity(t *testing.T) {
	// 13_loop_with_answer moved to testdata/examples/type-error (2026-08-22):
	// its no-declared-return function returns int (loop) and string (tail) —
	// the new cross-exit return-type consistency check (multi-return exits
	// are legal, value types must match) now rejects it.
	loopCases := []string{
		"09_iteration.ncoda",
		"12_loop_with_break.ncoda",
		"20_array_iteration_formatting.ncoda",
		"21_iteration_flatten_output.ncoda",
		"37_iteration_flatten_disabled.ncoda",
		"45_iteration_yield.ncoda",
		"46_loop_escape.ncoda",
		"49_for_statement_plain.ncoda",
		"for_expression.ncoda",
	}
	corpus := "../../compiler/testdata/examples/nodecoda"
	for _, name := range loopCases {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(corpus, name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			res := passes.DefaultPipeline().Run(string(src), name, nil)
			if res.HasErrors() || res.Program == nil {
				t.Fatalf("frontend rejected loop fixture")
			}
			graph, err := ir.Build(res.Program)
			if err != nil {
				t.Fatalf("IR build: %v", err)
			}
			gotAST, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
			if err != nil {
				t.Fatalf("IR render: %v", err)
			}
			if got, want := serialize(t, gotAST), readGolden(t, name+".yml"); got != want {
				t.Fatalf("IR loop render differs from frozen legacy golden")
			}
		})
	}
}

func TestIRRenderNonLoopParity(t *testing.T) {
	cases := []string{
		"04_code_execution.ncoda",
		"10_complex_multi_step.ncoda",
		"11_dual_switch_variable_aggregator.ncoda",
		"18_file_upload.ncoda",
		"19_http_request_json_tool.ncoda",
		"28_file_chat.ncoda",
		"29_ws2_workflow.ncoda",
		"57_parameter_extractor.ncoda",
		"customer-support.ncoda",
		"parallel_for_keep_null.ncoda",
	}
	corpus := "../../compiler/testdata/examples/nodecoda"
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(corpus, name))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			res := passes.DefaultPipeline().Run(string(src), name, nil)
			if res.HasErrors() || res.Program == nil {
				t.Fatalf("frontend rejected parity fixture")
			}
			graph, err := ir.Build(res.Program)
			if err != nil {
				t.Fatalf("IR build: %v", err)
			}
			gotAST, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
			if err != nil {
				t.Fatalf("IR render: %v", err)
			}
			if got, want := serialize(t, gotAST), readGolden(t, name+".yml"); got != want {
				t.Fatalf("IR non-loop render differs from frozen legacy golden")
			}
		})
	}
}

func TestIRRenderAttemptOperationParity(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kind string
	}{
		{
			name: "llm",
			kind: "llm",
			src: `function main() {
    attempt llm("openai_api_compatible/gpt-5.4", {"messages": [{"role": "user", "content": "hi"}]}) with retry(max: 2, interval: 1s) as result {
        success { return "ok"; }
        failure (err) { return "failed"; }
    }
}`,
		},
		{
			name: "tool",
			kind: "tool",
			src: `import "dify.time";
function main() {
    attempt time.current_time({"timezone": "UTC"}) with retry(max: 2, interval: 1s) as result {
        success { return "ok"; }
        failure (err) { return "failed"; }
    }
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res *passes.FrontendResult
			if tc.kind == "tool" {
				res = passes.DefaultPipeline().WithToolRegistry(timeCurrentTimeRegistry(t)).Run(tc.src, tc.name+"_attempt.ncoda", nil)
			} else {
				res = passes.DefaultPipeline().Run(tc.src, tc.name+"_attempt.ncoda", nil)
			}
			if res.HasErrors() || res.Program == nil {
				t.Fatalf("frontend rejected attempt: %v", res.Diagnostics)
			}
			graph, err := ir.Build(res.Program)
			if err != nil {
				t.Fatalf("IR build: %v", err)
			}
			attempt, ok := graph.Root.Stmts[0].(*ir.AttemptStmt)
			if !ok {
				t.Fatalf("root stmt = %T, want *ir.AttemptStmt", graph.Root.Stmts[0])
			}
			switch op := attempt.Op.(type) {
			case *ir.LLMCall:
				if op.Exception == nil || !op.Exception.FailBranch {
					t.Fatalf("LLM attempt lost failure branch: %#v", op.Exception)
				}
			case *ir.ToolCall:
				if op.Exception == nil || !op.Exception.FailBranch {
					t.Fatalf("tool attempt lost failure branch: %#v", op.Exception)
				}
			default:
				t.Fatalf("attempt operation = %T", attempt.Op)
			}

			got, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
			if err != nil {
				t.Fatalf("IR render: %v", err)
			}
			// llm attempt has a frozen legacy golden; the qualified tool
			// attempt (time.current_time) has no legacy snapshot (the
			// oracle cannot dispatch qualified calls) — the IR-level and
			// render-level assertions above lock the node instead.
			if tc.kind == "llm" {
				if gotYAML, wantYAML := serialize(t, got), readGolden(t, "attempt_llm.yml"); gotYAML != wantYAML {
					t.Fatalf("attempt llm render differs from frozen legacy golden")
				}
			}
			var strategy *string
			var retry *dify.RetryConfig
			for _, node := range got.Graph.Nodes {
				switch data := node.Data.(type) {
				case *dify.LLMDataNode:
					if tc.kind == "llm" {
						strategy = data.ErrorStrategy
						retry = data.RetryConfig
					}
				case *dify.ToolDataNode:
					if tc.kind == "tool" {
						strategy = data.ErrorStrategy
						retry = data.RetryConfig
					}
				}
			}
			if strategy == nil || *strategy != "fail-branch" {
				t.Fatalf("%s error strategy = %v, want fail-branch", tc.kind, strategy)
			}
			if retry == nil || retry.MaxRetries != 2 || retry.RetryInterval != 1000 || !retry.RetryEnabled {
				t.Fatalf("%s retry config = %#v, want 2 attempts/1000ms/enabled", tc.kind, retry)
			}
		})
	}
}

func TestIRRenderToolRetryParity(t *testing.T) {
	// Migrated to the qualified direct-call form in Task 4 (time is an
	// imported namespace + capability registry). The legacy lowering oracle
	// cannot dispatch qualified calls, so no golden baseline exists; the IR
	// retry envelope and the rendered RetryConfig below lock the node
	// (same assertions the legacy compare used to back, now IR-native).
	src := `import "dify.time";
function main() {
    let result = time.current_time({"timezone": "UTC"}) with retry(max: 2, interval: 1s);
    return "ok";
}`
	res := passes.DefaultPipeline().WithToolRegistry(timeCurrentTimeRegistry(t)).Run(src, "tool_retry.ncoda", nil)
	if res.HasErrors() || res.Program == nil {
		t.Fatalf("frontend rejected tool retry: %v", res.Diagnostics)
	}
	graph, err := ir.Build(res.Program)
	if err != nil {
		t.Fatalf("IR build: %v", err)
	}
	stmt, ok := graph.Root.Stmts[0].(*ir.NodeStmt)
	if !ok {
		t.Fatalf("root stmt = %T, want *ir.NodeStmt", graph.Root.Stmts[0])
	}
	tool, ok := stmt.Node.(*ir.ToolCall)
	if !ok || tool.Exception == nil || tool.Exception.MaxRetry != 2 || tool.Exception.RetryIntervalMS != 1000 {
		t.Fatalf("tool retry IR = %#v", stmt.Node)
	}

	got, err := dify.RenderFromIR(graph, res.Program.Program.Mode())
	if err != nil {
		t.Fatalf("IR render: %v", err)
	}
	var retry *dify.RetryConfig
	for _, node := range got.Graph.Nodes {
		if data, ok := node.Data.(*dify.ToolDataNode); ok {
			retry = data.RetryConfig
			if data.ToolName != "time" || data.ProviderID != "time" {
				t.Fatalf("tool node identity = %q/%q, want time/time", data.ProviderID, data.ToolName)
			}
		}
	}
	if retry == nil || retry.MaxRetries != 2 || retry.RetryInterval != 1000 || !retry.RetryEnabled {
		t.Fatalf("rendered retry config = %#v, want 2 attempts/1000ms/enabled", retry)
	}
}

// timeCurrentTimeRegistry is the closed-world capability definition behind
// the qualified time.current_time fixtures (mirrors the Task 4 capability
// header internal/compiler/testdata/examples/capabilities/time.ncoda, which
// declares retry+failure_branch support).
func timeCurrentTimeRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	registry, err := tool.NewRegistry(tool.Definition{
		Provider:              "time",
		Operation:             "current_time",
		Parameters:            []tool.Parameter{{Name: "timezone", Type: "any"}},
		SupportsRetry:         true,
		SupportsFailureBranch: true,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func serialize(t *testing.T, ast *dify.WorkflowAST) string {
	t.Helper()
	om, err := dify.SerializeToDict(ast)
	if err != nil {
		t.Fatalf("SerializeToDict: %v", err)
	}
	yml, err := dify.SerializeYAML(om)
	if err != nil {
		t.Fatalf("SerializeYAML: %v", err)
	}
	return yml
}
