package trace

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestFileWriterEnrichesAndPersists(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileWriter(dir, Meta{Workflow: "flow_1", Profile: "placeholder", SemanticsVersion: "v1"})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	rec := Record{Stage: "fill", RuleID: RuleFill, Origin: OriginPolicy, Evidence: "hand:docs/x.md:1", FillRule: "absent_scalar_zero", Attempt: 1, Axis: "total", Input: "x", Output: "filled", Decision: "filled"}
	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := filepath.Base(w.Path()); got != "flow_1.jsonl" {
		t.Fatalf("path = %q, want flow_1.jsonl", got)
	}
	f, err := os.Open(w.Path())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatal("no record written")
	}
	var got Record
	if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Workflow != "flow_1" || got.Profile != "placeholder" || got.SemanticsVersion != "v1" {
		t.Fatalf("metadata not stamped: %+v", got)
	}
	if err := got.ValidateFull(); err != nil {
		t.Fatalf("full record: %v", err)
	}
}

func TestFileWriterConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileWriter(dir, Meta{Workflow: "conc"})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Write(Record{Stage: "retry", RuleID: RuleRetry, Origin: OriginWireAbsent, Evidence: "hand:docs/x.md:1", Attempt: 1, Axis: "total"})
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := 0
	for _, b := range raw {
		if b == '\n' {
			lines++
		}
	}
	if lines != 50 {
		t.Fatalf("lines = %d, want 50", lines)
	}
}

func TestValidateFullRejectsEachMissingField(t *testing.T) {
	base := Record{Stage: "timeout", RuleID: RuleTimeout, Origin: OriginWireAbsent, Evidence: "hand:docs/x.md:1", FillRule: "timeout_axis", Attempt: 1, Axis: "total", Input: "in", Output: "out"}
	if err := base.ValidateFull(); err != nil {
		t.Fatalf("base should be full: %v", err)
	}
	cases := []struct {
		name string
		drop func(*Record)
	}{
		{"fill_rule", func(r *Record) { r.FillRule = "" }},
		{"attempt", func(r *Record) { r.Attempt = 0 }},
		{"axis", func(r *Record) { r.Axis = "" }},
		{"input", func(r *Record) { r.Input = "" }},
		{"output", func(r *Record) { r.Output = "" }},
	}
	for _, tc := range cases {
		r := base
		tc.drop(&r)
		if err := r.ValidateFull(); err == nil {
			t.Fatalf("dropping %s still validated", tc.name)
		}
	}
}

func TestFileWriterSourceKeyKeepsSameStemApart(t *testing.T) {
	dir := t.TempDir()
	w1, err := NewFileWriter(dir, Meta{Workflow: "flow", SourceKey: "/corpus/a/flow.spec.json"})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer w1.Close()
	w2, err := NewFileWriter(dir, Meta{Workflow: "flow", SourceKey: "/corpus/b/flow.spec.json"})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer w2.Close()
	if w1.Path() == w2.Path() {
		t.Fatalf("same stem and different sources share one trace file: %s", w1.Path())
	}
	if got := filepath.Base(w1.Path()); got == "flow.jsonl" || !strings.HasPrefix(got, "flow__") || !strings.HasSuffix(got, ".jsonl") {
		t.Fatalf("hashed trace name = %q", got)
	}
	if err := w1.Write(Record{Stage: "load", RuleID: RuleFill, Origin: OriginPolicy, Evidence: "hand:docs/x.md:1", FillRule: "r", Attempt: 1, Axis: "total"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Same source must be stable across writers (re-runs overwrite one file).
	w3, err := NewFileWriter(dir, Meta{Workflow: "flow", SourceKey: "/corpus/a/flow.spec.json"})
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	defer w3.Close()
	if w3.Path() != w1.Path() {
		t.Fatalf("same source produced different paths: %s != %s", w3.Path(), w1.Path())
	}
}

func TestFileWriterRefusesOutOfRootSymlink(t *testing.T) {
	dir := t.TempDir()
	meta := Meta{Workflow: "flow", SourceKey: "/corpus/a/flow.spec.json"}
	w, err := NewFileWriter(dir, meta)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	path := w.Path()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	victim := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatalf("victim: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove trace: %v", err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if w2, err := NewFileWriter(dir, meta); err == nil {
		_ = w2.Close()
		t.Fatal("writer followed a symlink out of the trace dir")
	}
	raw, err := os.ReadFile(victim)
	if err != nil || string(raw) != "keep" {
		t.Fatalf("victim clobbered: %q (err %v)", raw, err)
	}
}
