package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/nodecoda/ncoda/pkg/migrate"
)

const cliSource = `@language nodecoda/1
type Answer = { string answer; };
function main() -> string { let r = llm<Answer>("m", {}); return r.answer; }
`

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunDefaultAndCheckNeverWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.ncoda")
	writeTestFile(t, path, cliSource, 0640)

	code, stdout, stderr := invoke(t, path)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("default = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	data, _ := os.ReadFile(path)
	if string(data) != cliSource {
		t.Fatal("default mode wrote source")
	}

	code, stdout, stderr = invoke(t, "--check", path)
	if code != 1 || stdout != "" || stderr != "" {
		t.Fatalf("check = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	data, _ = os.ReadFile(path)
	if string(data) != cliSource {
		t.Fatal("check mode wrote source")
	}
}

func TestRunStdoutAndUsageValidation(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ncoda")
	b := filepath.Join(dir, "b.ncoda")
	writeTestFile(t, a, cliSource, 0644)
	writeTestFile(t, b, cliSource, 0644)

	code, stdout, stderr := invoke(t, "--stdout", a)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "r.text.answer") {
		t.Fatalf("stdout = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, args := range [][]string{
		{"--stdout", a, b},
		{"--stdout", "--in-place", a},
		{"--diagnostics", "xml", a},
	} {
		code, _, _ = invoke(t, args...)
		if code != 2 {
			t.Fatalf("args %v: code %d, want 2", args, code)
		}
	}
}

func TestRunInPlaceDryRunAndModePreservation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workflow.ncoda")
	writeTestFile(t, path, cliSource, 0750)

	code, _, stderr := invoke(t, "--in-place", "--dry-run", path)
	if code != 0 || stderr != "" {
		t.Fatalf("dry run = code %d stderr %q", code, stderr)
	}
	data, _ := os.ReadFile(path)
	if string(data) != cliSource {
		t.Fatal("dry-run wrote source")
	}

	code, _, stderr = invoke(t, "--in-place", path)
	if code != 0 || stderr != "" {
		t.Fatalf("in-place = code %d stderr %q", code, stderr)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "r.text.answer") {
		t.Fatalf("in-place did not rewrite:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0750 {
		t.Fatalf("mode = %o, want 750", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary sibling leaked: %v", entries)
	}
}

func TestRunBatchInnerMarkerIsProductionProvenance(t *testing.T) {
	dir := t.TempDir()
	tagged := filepath.Join(dir, "tagged.ncoda")
	untagged := filepath.Join(dir, "untagged.ncoda")
	taggedSource := `@language nodecoda/1
type Answer = { string $output; };
function main() -> string {
    let renamed_batch_value = ` + migrate.BatchInnerSourceMarker + ` llm<Answer>("m", {});
    return renamed_batch_value.$output;
}
`
	untaggedSource := strings.ReplaceAll(taggedSource,
		"renamed_batch_value = "+migrate.BatchInnerSourceMarker,
		"ordinary_inner =")
	untaggedSource = strings.ReplaceAll(untaggedSource, "renamed_batch_value.$output", "ordinary_inner.$output")
	writeTestFile(t, tagged, taggedSource, 0o644)
	writeTestFile(t, untagged, untaggedSource, 0o644)

	code, stdout, stderr := invoke(t, "--stdout", tagged)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "renamed_batch_value.text") {
		t.Fatalf("tagged migration = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke(t, "--stdout", untagged)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ordinary_inner.$output") || strings.Contains(stdout, "ordinary_inner.text") {
		t.Fatalf("untagged migration = code %d stdout %q stderr %q", code, stdout, stderr)
	}
}

func TestRunRecursiveOrderingAndJSONDiagnostics(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "a.ncoda")
	b := filepath.Join(dir, "b.ncoda")
	bad := `@language nodecoda/1
type Answer = { string answer; };
function main(bool choose) -> string {
 let a = llm<Answer>("m", {}); let b = llm<Answer>("m", {}); var r;
 if (choose) { r = a; } else { r = b; }
 return r.answer;
}`
	writeTestFile(t, b, bad, 0644)
	writeTestFile(t, a, bad, 0644)
	writeTestFile(t, filepath.Join(dir, "ignored.txt"), bad, 0644)

	code, stdout, stderr := invoke(t, "--diagnostics", "json", dir)
	if code != 1 || stderr != "" {
		t.Fatalf("json diagnostics = code %d stdout %q stderr %q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSON lines = %d: %q", len(lines), stdout)
	}
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		for _, key := range []string{"path", "line", "column", "code", "severity", "message"} {
			if _, ok := item[key]; !ok {
				t.Fatalf("missing %s in %v", key, item)
			}
		}
		paths = append(paths, item["path"].(string))
	}
	if !sort.StringsAreSorted(paths) || paths[0] != a || paths[1] != b {
		t.Fatalf("paths not lexical: %v", paths)
	}
}

func TestRunTextDiagnosticAndNoPartialRewrite(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.ncoda")
	bad := filepath.Join(dir, "bad.ncoda")
	badSource := "function main( {"
	writeTestFile(t, good, cliSource, 0644)
	writeTestFile(t, bad, badSource, 0600)

	code, _, stderr := invoke(t, "--in-place", good, bad)
	if code != 1 || !strings.Contains(stderr, "bad.ncoda:1:") || !strings.Contains(stderr, "LLM_MIGRATE_PARSE") {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
	badData, _ := os.ReadFile(bad)
	if string(badData) != badSource {
		t.Fatal("failed file was partially rewritten")
	}
	goodData, _ := os.ReadFile(good)
	if !strings.Contains(string(goodData), "r.text.answer") {
		t.Fatal("independent good file was not rewritten")
	}
}

func TestRunIOFailureIsExitTwo(t *testing.T) {
	code, _, stderr := invoke(t, filepath.Join(t.TempDir(), "missing.ncoda"))
	if code != 2 || stderr == "" {
		t.Fatalf("code %d stderr %q", code, stderr)
	}
}
