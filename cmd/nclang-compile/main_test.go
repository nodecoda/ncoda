package main

// main_test.go — the language-lane CLI's contract smoke.
//
// The open distribution ships the language lane only (ASSET-BOUNDARY P2c):
// stdin source → ncoda-ir/v1 on stdout. Exit codes: 0 ok · 1 language red ·
// 2 usage (including any platform target).

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/irdump"
)

func runCLITest(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	_ = errOut
	code := runCLI(strings.NewReader(stdin), &out, args)
	return code, out.String(), errOut.String()
}

func TestLanguageBuildVersion(t *testing.T) {
	code, out, _ := runCLITest(t, "", "--version")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.HasPrefix(out, "nclang-compile ") {
		t.Fatalf("version output = %q", out)
	}
	if !strings.Contains(out, "(contract ") {
		t.Fatalf("version output must carry the contract version: %q", out)
	}
}

func TestLanguageBuildEmptyInputIsRed(t *testing.T) {
	code, out, _ := runCLITest(t, "")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (language red)", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("a rejected program must not print a dump: %q", out)
	}
}

func TestLanguageBuildMinimalProgramEmitsIR(t *testing.T) {
	src := "@language nodecoda/1\n@mode workflow\nfunction main() {\n    output(\"hello\");\n}\n"
	code, out, _ := runCLITest(t, src)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	var dump map[string]any
	if err := json.Unmarshal([]byte(out), &dump); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if dump["schema"] != irdump.IRDumpSchema {
		t.Fatalf("schema = %v, want %s", dump["schema"], irdump.IRDumpSchema)
	}
}

func TestLanguageBuildPlatformTargetIsUsageError(t *testing.T) {
	code, _, _ := runCLITest(t, "", "--target", "dify-1.16-graphon-0.6")
	if code != 2 {
		t.Fatalf("exit=%d, want 2: the open distribution ships no platform adapter", code)
	}
}

func TestLanguageBuildUnknownFlagIsUsageError(t *testing.T) {
	code, _, _ := runCLITest(t, "", "--coze-biz-out", "x")
	if code != 2 {
		t.Fatalf("exit=%d, want 2 for an undefined flag", code)
	}
}

func TestLanguageBuildContractShapeWithoutIR(t *testing.T) {
	src := "@language nodecoda/1\n@mode workflow\nfunction main() {\n    output(\"hello\");\n}\n"
	code, out, _ := runCLITest(t, src, "--emit-ir=false")
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got["status"] != "ok" || got["ok"] != true {
		t.Fatalf("status=%v ok=%v, want ok/true", got["status"], got["ok"])
	}
	if _, hasIR := got["ir"]; hasIR {
		t.Fatal("--emit-ir=false must not carry an IR dump")
	}
}
