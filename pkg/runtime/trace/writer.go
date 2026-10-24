package trace

// writer.go — production trace sink (P5).
//
// The trace package owns the record shape and validation; this file owns the
// only on-disk sink used by `ncoda run` (ES-062/ES-064). Each workflow run gets
// one JSONL file named from the artifact stem, so concurrent 96-case runs never
// interleave records. The writer enriches every record with the run metadata
// (workflow/profile/semantics_version) that nodes cannot know, and it never
// rewrites a field a node already set.
//
// Redaction boundary: nodes only put port names, rule ids, decisions and
// numeric budgets into Input/Output (see the emit* call sites). The writer does
// not accept payload values, so a fixture URL/key/user field cannot reach disk
// through this path. The P5 gate still scans the produced files for markers.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// RuleHarness records that the run is using a declared harness profile
// (echo model / stub tools / placeholder inputs). It is the machine-readable
// half of ES-061: a harness value must never masquerade as platform evidence.
const RuleHarness = "SEM-HARNESS-001"

// HarnessEvidence is the resolvable pointer to the declared deviations file.
const HarnessEvidence = "hand:docs/five-step/exec-semantics/harness-profile.yaml:1"

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// Meta is the per-run metadata stamped onto every record.
//
// SourceKey is the run's identity (the artifact path). When set, the file name
// carries a short hash of it, so two artifacts that share a base name in
// different directories cannot silently overwrite each other's trace.
type Meta struct {
	Workflow         string
	Profile          string
	SemanticsVersion string
	SourceKey        string
}

// FileWriter appends validated-shape trace records to one JSONL file.
type FileWriter struct {
	mu   sync.Mutex
	f    *os.File
	root *os.Root
	meta Meta
	path string
	err  error
}

// NewFileWriter creates <dir>/<workflow>.jsonl and returns the sink. workflow
// is sanitized to a single path component; empty becomes "workflow".
func NewFileWriter(dir string, meta Meta) (*FileWriter, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("trace: empty trace dir")
	}
	name := strings.TrimSpace(meta.Workflow)
	if name == "" {
		name = "workflow"
	}
	name = unsafeFileChars.ReplaceAllString(name, "_")
	if name == "" || name == "." || name == ".." {
		name = "workflow"
	}
	if key := strings.TrimSpace(meta.SourceKey); key != "" {
		sum := sha256.Sum256([]byte(key))
		name += "__" + hex.EncodeToString(sum[:4])
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("trace: create dir: %w", err)
	}
	path := filepath.Join(dir, name+".jsonl")
	// Open through an os.Root: the sink only ever resolves a single path
	// component inside the trace dir, so a pre-planted symlink in a shared
	// temp directory cannot redirect the write.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("trace: open dir: %w", err)
	}
	f, err := root.OpenFile(name+".jsonl", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("trace: open %s: %w", path, err)
	}
	return &FileWriter{f: f, root: root, meta: meta, path: path}, nil
}

// Path returns the on-disk trace path.
func (w *FileWriter) Path() string { return w.path }

// Write enriches and appends one record. Write is safe for concurrent node
// callbacks. It never mutates the caller's record.
func (w *FileWriter) Write(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	if w.f == nil {
		w.err = fmt.Errorf("trace: writer is closed")
		return w.err
	}
	if r.Workflow == "" {
		r.Workflow = w.meta.Workflow
	}
	if r.Profile == "" {
		r.Profile = w.meta.Profile
	}
	if r.SemanticsVersion == "" {
		r.SemanticsVersion = w.meta.SemanticsVersion
	}
	line, err := json.Marshal(r)
	if err != nil {
		w.err = fmt.Errorf("trace: marshal: %w", err)
		return w.err
	}
	if _, err := w.f.Write(append(line, '\n')); err != nil {
		w.err = fmt.Errorf("trace: write: %w", err)
		return w.err
	}
	return nil
}

// Close flushes and closes the file. It returns the first write error so the
// CLI can warn without changing the run's exit code (trace is observation).
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil && w.root == nil {
		return w.err
	}
	if w.f != nil {
		if err := w.f.Close(); err != nil && w.err == nil {
			w.err = err
		}
		w.f = nil
	}
	if w.root != nil {
		if err := w.root.Close(); err != nil && w.err == nil {
			w.err = err
		}
		w.root = nil
	}
	return w.err
}
