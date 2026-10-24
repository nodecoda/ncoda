package codeparse

import (
	"strings"
	"testing"
)

func TestCheckPythonSyntax(t *testing.T) {
	ok, vs, err := CheckOne(Item{ID: "n1", Lang: "python",
		Code: "async def main(args):\n    return {\"x\": 1}\n"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !ok.OK {
		t.Fatalf("valid python reported as error: %s", ok.Err)
	}
	if vs.Python == "" {
		t.Fatal("python version not observed")
	}

	bad, _, err := CheckOne(Item{ID: "n2", Lang: "python",
		Code: "async def main(args) -> dict:\n    return {\"a\": 1 b}\n"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if bad.OK {
		t.Fatal("invalid python reported as ok")
	}
	if !strings.Contains(bad.Err, "SyntaxError") {
		t.Fatalf("error = %q, want SyntaxError", bad.Err)
	}
}

// TestCheckNeverExecutes is the safety boundary: compile-only must not run
// user code. Both snippets would be observable (exit / infinite loop) if the
// checker executed them.
func TestCheckNeverExecutes(t *testing.T) {
	for _, code := range []string{
		"async def main(args):\n    raise SystemExit(3)\n",
		"async def main(args):\n    while True:\n        pass\n",
	} {
		r, _, err := CheckOne(Item{ID: "n", Lang: "python", Code: code})
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if !r.OK {
			t.Fatalf("compile-only rejected valid source: %s", r.Err)
		}
	}
}

func TestCheckJSSyntaxWithPlatformSignature(t *testing.T) {
	// The platform template is TypeScript-shaped; the checker must apply the same
	// strip rule as the executor, or every node would be a false positive.
	ok, vs, err := CheckOne(Item{ID: "j1", Lang: "javascript",
		Code: "async function main({ params }: Args): Promise<Output> {\n  return { a: 1 };\n}\n"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !ok.OK {
		t.Fatalf("valid platform JS rejected: %s", ok.Err)
	}
	if vs.Node == "" {
		t.Fatal("node version not observed")
	}

	bad, _, err := CheckOne(Item{ID: "j2", Lang: "javascript",
		Code: "async def main(args):\n  return {}\n"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if bad.OK {
		t.Fatal("python-in-js reported as ok")
	}
}

// TestCheckJSAcceptsESMAndRejectsTopLevelReturn locks the two goals the
// runtime actually accepts: `node -` auto-detects ESM (import/export), while
// a bare top-level `return` fails under both the script and module goals.
func TestCheckJSAcceptsESMAndRejectsTopLevelReturn(t *testing.T) {
	esm, _, err := CheckOne(Item{ID: "j3", Lang: "javascript",
		Code: "import pad from 'leftpad';\nasync function main({ params }) { return { out: pad(params.input) }; }\n"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !esm.OK {
		t.Fatalf("valid ESM source rejected: %s", esm.Err)
	}

	bare, _, err := CheckOne(Item{ID: "j4", Lang: "javascript",
		Code: "return { a: 1 };\n"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if bare.OK {
		t.Fatal("top-level return is not runnable by the node executor; must be red")
	}
	if !strings.Contains(strings.ToLower(bare.Err), "return statement is not allowed") {
		t.Fatalf("error = %q, want a top-level-return rejection", bare.Err)
	}
}

func TestCheckBatchGroupsLanguages(t *testing.T) {
	items := []Item{
		{ID: "a", Lang: "python", Code: "x = 1\n"},
		{ID: "b", Lang: "javascript", Code: "const x = 1;\n"},
		{ID: "c", Lang: "python", Code: "def f(:\n"},
	}
	rs, vs, err := Check(items)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(rs) != 3 {
		t.Fatalf("results = %d, want 3", len(rs))
	}
	got := map[string]bool{}
	for _, r := range rs {
		got[r.ID] = r.OK
	}
	if !got["a"] || !got["b"] || got["c"] {
		t.Fatalf("verdicts = %v, want a,b ok and c red", got)
	}
	if vs.Python == "" || vs.Node == "" {
		t.Fatalf("versions incomplete: %+v", vs)
	}
}

func TestBlacklistedPythonModules(t *testing.T) {
	if hits := BlacklistedPythonModules("import json\nimport socket\n"); len(hits) != 1 || hits[0] != "socket" {
		t.Fatalf("hits = %v, want [socket]", hits)
	}
	if hits := BlacklistedPythonModules("# import socket\nimport json\n"); len(hits) != 0 {
		t.Fatalf("comment import counted: %v", hits)
	}
	if hits := BlacklistedPythonModules("from threading import Thread\n"); len(hits) != 1 || hits[0] != "threading" {
		t.Fatalf("hits = %v, want [threading]", hits)
	}
}

func TestSameMajorMinor(t *testing.T) {
	if SameMajorMinor("3.12.3", "3.9") {
		t.Fatal("3.12 vs 3.9 must differ in major.minor")
	}
	if !SameMajorMinor("3.9.18", "3.9") {
		t.Fatal("3.9.18 vs 3.9 must match")
	}
}
