package main

// main_dify_test.go — 开放发行版（-tags dify）二进制的契约冒烟。
// 闭源全量 CLI 的测试面在 main_test.go（!dify）；这里只锁开放面：
// 版本输出、空输入的 COMPILE_ERROR、最小程序的 SUCCEEDED、未知 flag 的用法错。

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runDifyCLI(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := runCLI(strings.NewReader(stdin), &out, args)
	return code, out.String()
}

func TestDifyBuildVersion(t *testing.T) {
	code, out := runDifyCLI(t, "", "--version")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.HasPrefix(out, "nclang-compile ") {
		t.Fatalf("version output = %q", out)
	}
}

func TestDifyBuildEmptyInputIsCompileError(t *testing.T) {
	code, out := runDifyCLI(t, "")
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (contract results use exit 0)", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got["status"] != "COMPILE_ERROR" {
		t.Fatalf("status = %v, want COMPILE_ERROR", got["status"])
	}
}

func TestDifyBuildMinimalProgramSucceeds(t *testing.T) {
	src := "@language nodecoda/1\n@mode workflow\nfunction main() {\n    output(\"hello\");\n}\n"
	code, out := runDifyCLI(t, src)
	if code != 0 {
		t.Fatalf("exit=%d, want 0: %s", code, out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got["status"] != "SUCCEEDED" || got["ok"] != true {
		t.Fatalf("status=%v ok=%v, want SUCCEEDED/true", got["status"], got["ok"])
	}
	if y, _ := got["yaml"].(string); y == "" {
		t.Fatalf("yaml is empty: %s", out)
	}
}

func TestDifyBuildUnknownFlagIsUsageError(t *testing.T) {
	code, _ := runDifyCLI(t, "", "--coze-biz-out", "x")
	if code != 2 {
		t.Fatalf("exit=%d, want 2 for an undefined flag", code)
	}
}
