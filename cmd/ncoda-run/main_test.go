package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rc contract is the CLI's public surface: 0 result, 1 language/run
// failure, 2 usage. These tests pin all three against the real entry point.
func TestRunCLIContract(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		args   []string
		wantRC int
		want   string
	}{
		{
			name:   "minimal source runs end to end",
			src:    "function main() -> string { return \"hi\"; }",
			wantRC: 0,
			want:   `"output":"hi"`,
		},
		{
			name:   "syntax error is a language failure",
			src:    "function main( {",
			wantRC: 1,
			want:   "SYNTAX_ERROR",
		},
		{
			name:   "unknown model factory is a usage error",
			src:    "function main() -> string { return \"hi\"; }",
			args:   []string{"--model-factory", "openai"},
			wantRC: 2,
			want:   "not available in the open distribution",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			rc := runCLI(strings.NewReader(c.src), &out, &errb, c.args)
			if rc != c.wantRC {
				t.Fatalf("rc = %d, want %d (stderr: %s)", rc, c.wantRC, errb.String())
			}
			got := out.String() + errb.String()
			if !strings.Contains(got, c.want) {
				t.Fatalf("output %q missing %q", got, c.want)
			}
		})
	}
}

func TestRunCLIInputAndSources(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "hello.ncoda")
	if err := os.WriteFile(src, []byte("function main(string who) -> string { return `hi ${who}`; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if rc := runCLI(nil, &out, &errb, []string{src, "-p", "who=ada", "--raw"}); rc != 0 {
		t.Fatalf("rc = %d, stderr=%s", rc, errb.String())
	}
	if got := strings.TrimSpace(out.String()); got != "hi ada" {
		t.Fatalf("raw output = %q, want %q", got, "hi ada")
	}

	specPath := filepath.Join(dir, "spec.json")
	out.Reset()
	if rc := runCLI(strings.NewReader("function main() -> string { return \"x\"; }"), &out, &errb, []string{"--emit-spec", specPath}); rc != 0 {
		t.Fatalf("emit-spec rc = %d, stderr=%s", rc, errb.String())
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("emit-spec is not JSON: %v", err)
	}
	if _, ok := doc["nodes"]; !ok {
		t.Fatalf("emit-spec missing nodes: %s", raw)
	}
}
