package dify_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/backend/dify"
)

// referencePython locates the CPython reference interpreter: the project
// venv first, then python3/python on PATH, honoring NCLANG_PYTHON as an
// explicit override. Tests skip when no interpreter is available.
func referencePython(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("NCLANG_PYTHON"); bin != "" {
		// The override must also point at a real interpreter: a mis-set
		// NCLANG_PYTHON skips (with the same message as the candidate path)
		// instead of failing later with a confusing exec error.
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("python reference unavailable: NCLANG_PYTHON=%q: %v", bin, err)
		}
		return bin
	}
	candidates := []string{"<reference-venv>/bin/python"}
	for _, name := range []string{"python3", "python"} {
		if bin, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, bin)
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Skip("python reference unavailable; set NCLANG_PYTHON to run parity tests")
	return ""
}

// jsonDumpsString must be byte-identical to CPython json.dumps(s,
// ensure_ascii=True) for a broad corpus (the code_materializer.py:84
// StringLit rendering). The corpus is shipped to Python as JSON so no
// source quoting can distort control/unicode bytes.
func TestJSONDumpsStringParity(t *testing.T) {
	corpus := []string{
		"hello", "it's", `a"b`, "a\\b", "a\nb", "\t\r\b\f", "\x01", "\x7f",
		"你好，世界", "😀", "\u2028", "\x85", "line1\nline2", "a/b c~", "e\u0301",
	}
	payload, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(referencePython(t), "-c",
		"import json,sys; [print(json.dumps(s, ensure_ascii=True)) for s in json.load(sys.stdin)]")
	cmd.Stdin = strings.NewReader(string(payload))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(corpus) {
		t.Fatalf("python lines %d != corpus %d", len(lines), len(corpus))
	}
	for i, s := range corpus {
		want := lines[i]
		got := dify.JSONDumpsString(s)
		if got != want {
			t.Errorf("JSONDumpsString(%q) = %q, want %q", s, got, want)
		}
	}
}
