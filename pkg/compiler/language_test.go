package compiler

// language_test.go — 语言 lane（`--target __none__` + `--emit-ir`）与 P6 投影。

import (
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/irdump"
)

const languageHeader = `@language nodecoda/1
@mode workflow

type R = { string a; };
`

func compileLanguage(t *testing.T, body string, emitIR bool) *Result {
	t.Helper()
	return Compile(languageHeader+body, Options{
		Target:   TargetLanguageOnly,
		Filename: "language.ncoda",
		EmitIR:   emitIR,
	})
}

func hasDiagCode(res *Result, code string) bool {
	for _, d := range res.Diagnostics {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestLanguageLaneEmitsIRDump(t *testing.T) {
	res := compileLanguage(t, "function main(string p) -> R {\n    return {\"a\": p};\n}\n", true)
	if !res.OK {
		t.Fatalf("language lane rejected a valid program: %+v", res.Diagnostics)
	}
	if res.Status != "ok" {
		t.Fatalf("status = %q, want ok (the language lane has no platform product)", res.Status)
	}
	if res.IR == nil {
		t.Fatal("--emit-ir produced no dump")
	}
	if res.IR.Schema != irdump.IRDumpSchema {
		t.Fatalf("schema = %q, want %q", res.IR.Schema, irdump.IRDumpSchema)
	}
	data, err := res.IR.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"schema": "ncoda-ir/v1"`) {
		t.Fatalf("dump does not carry the schema: %s", data)
	}
}

func TestLanguageLaneOmitsDumpUnlessRequested(t *testing.T) {
	res := compileLanguage(t, "function main() -> R {\n    return {\"a\": \"x\"};\n}\n", false)
	if !res.OK {
		t.Fatalf("language lane rejected a valid program: %+v", res.Diagnostics)
	}
	if res.IR != nil {
		t.Fatal("IR populated without EmitIR")
	}
}

func TestLanguageLaneRejectsPlatformTargets(t *testing.T) {
	// The open distribution has no platform backend: a platform target is
	// UNKNOWN_TARGET here (rc=2 usage), never a silent dify render.
	res := Compile(languageHeader+"function main() -> R {\n    return {\"a\": \"x\"};\n}\n",
		Options{Target: "coze-biz-1.0", Filename: "lang.ncoda"})
	if res.OK {
		t.Fatal("platform target accepted by the open distribution")
	}
	if res.Status != StatusUnknownTarget {
		t.Fatalf("status = %q, want %q", res.Status, StatusUnknownTarget)
	}
}

func TestCodeSyntaxProjectionIsALanguageFact(t *testing.T) {
	src := "function main() {\n" +
		"    let v = foreign code python3() -> R { source `async def main(args):\n" +
		"    ret = {\"a\": 1 b}\n" +
		"    return ret`; };\n" +
		"    return \"done\";\n}\n"
	res := compileLanguage(t, src, true)
	if res.OK {
		t.Fatal("invalid python source compiled OK in the language lane")
	}
	if !hasDiagCode(res, DiagCodeCodeSyntax) {
		t.Fatalf("missing CODE_SYNTAX: %+v", res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if d.Code == DiagCodeCodeSyntax && !strings.Contains(d.Message, "SyntaxError") {
			t.Fatalf("message = %q, want the parser error", d.Message)
		}
	}
}

func TestCodeSyntaxProjectionAcceptsValidSource(t *testing.T) {
	src := "function main() {\n" +
		"    let v = foreign code python3() -> R { source `def main() -> dict:\n" +
		"    return {\"a\": 1}`; };\n" +
		"    return \"done\";\n}\n"
	res := compileLanguage(t, src, true)
	if !res.OK {
		t.Fatalf("valid source rejected: %+v", res.Diagnostics)
	}
	if hasDiagCode(res, DiagCodeCodeSyntax) {
		t.Fatalf("CODE_SYNTAX on valid source: %+v", res.Diagnostics)
	}
}
